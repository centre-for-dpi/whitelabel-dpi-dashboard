package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/a11y"
)

// The structural accessibility gate.
//
// This runs the real binary's wiring through the same httptest harness as
// server_e2e_test.go and audits the HTML that comes out. It sits between two
// other layers: config validation checks the palette without seeing markup, and
// the browser suite under test/a11y checks the rendered result but needs Chrome.
// Everything decidable from the HTML alone belongs here, because this is the
// layer that runs on every commit.
//
// Every rule it applies was broken in this repository at least once. The
// aria-label-on-a-roleless-span rule, for instance, exists because the rank
// movement indicator carried its label that way in four rows of every locale,
// and no test noticed until axe-core was pointed at a running server.

// auditPath fetches a path and returns any structural problems in it.
func auditPath(t *testing.T, h http.Handler, path string) []a11y.Problem {
	t.Helper()
	body := get(t, h, path)
	problems, err := a11y.Audit(body)
	if err != nil {
		t.Fatalf("GET %s: parsing the response: %v", path, err)
	}
	return problems
}

// pagePaths are the full documents a reader can land on. Fragments are audited
// separately because they are not whole documents and the document-level rules
// do not apply to them.
func pagePaths() []string {
	return []string{
		"/",
		"/?scope=state",
		"/?theme=dark",
		"/?q=pension",
		"/?q=zzzznothingmatches",     // the empty state
		"/?status=major,partial",     // filtered
		"/?sort=name&dir=desc",       // sorted
		"/service/pan",               // drawer open, full page
		"/service/pan?tab=errors",    // a tab with a disclosure in it
		"/service/pan?tab=traffic",   // a tab with a chart and a table
		"/service/pan?tab=incidents", // a tab with a timeline
	}
}

func TestEveryPageIsStructurallyAccessible(t *testing.T) {
	h := dashboard(t)

	for _, path := range pagePaths() {
		t.Run(path, func(t *testing.T) {
			for _, p := range auditPath(t, h, path) {
				t.Errorf("%s", p)
			}
		})
	}
}

// Locale matters structurally, not just textually: a term that resolves in
// English and not in Arabic can empty an accessible name, and a right-to-left
// document must still declare its direction.
func TestEveryLocaleIsStructurallyAccessible(t *testing.T) {
	h := dashboard(t)

	for _, locale := range []string{"en", "ar", "hi", "zh", "es", "fr", "ru", "sw"} {
		t.Run(locale, func(t *testing.T) {
			for _, path := range []string{"/", "/service/pan"} {
				for _, p := range auditPath(t, h, path+"?lang="+locale) {
					t.Errorf("%s%s: %s", path, "?lang="+locale, p)
				}
			}
		})
	}
}

// Fragments are swapped into a live document, so their own markup has to hold up
// even though they are not documents themselves. The document-level rules —
// title, lang — cannot apply, so they are filtered out rather than asserted.
func TestEveryFragmentIsStructurallyAccessible(t *testing.T) {
	h := dashboard(t)

	documentOnly := map[string]bool{
		"document-title": true,
		"html-lang":      true,
		"html-dir":       true,
		"heading-h1":     true,
	}

	for _, path := range []string{
		"/fragments/leaderboard",
		"/fragments/leaderboard?q=pension",
		"/fragments/leaderboard?q=zzzznothingmatches",
		"/fragments/verdict",
		"/fragments/signals",
		"/fragments/service/pan",
		"/fragments/service/pan?tab=errors",
		"/fragments/service/pan?tab=traffic",
		"/fragments/service/pan?tab=incidents",
	} {
		t.Run(path, func(t *testing.T) {
			for _, p := range auditPath(t, h, path) {
				if documentOnly[p.Rule] {
					continue
				}
				t.Errorf("%s", p)
			}
		})
	}
}

// A term id that resolves to itself renders the id on screen. The resolver does
// that deliberately — literal text in a config file simply works — but it means
// a typo is displayed rather than reported, and "flt.search" is not a label.
//
// server_e2e_test.go has a version of this check whose regex requires the id to
// sit immediately after '>', so it missed ids inside attributes and ids preceded
// by whitespace. footer.cdpi rendered in the footer of every locale for exactly
// that reason. This one looks at text and attribute values alike.
func TestNoTermIDReachesTheReader(t *testing.T) {
	h := dashboard(t)

	// Prefixes that are term-id namespaces in this repository's locale files.
	prefixes := []string{
		"brand.", "chrome.", "verdict.", "legend.", "sig.", "lb.", "flt.",
		"dr.", "metric.", "status.", "rule.", "period.", "inc.", "footer.",
		"theme.", "cat.", "req.",
	}

	for _, locale := range []string{"en", "ar", "hi", "zh", "es", "fr", "ru", "sw"} {
		for _, path := range []string{"/", "/service/pan", "/service/pan?tab=incidents"} {
			url := path + ampOrQ(path) + "lang=" + locale
			leaks, err := a11y.TermIDLeaks(get(t, h, url), prefixes)
			if err != nil {
				t.Fatalf("GET %s: %v", url, err)
			}
			for _, leak := range leaks {
				t.Errorf("%s renders the term id %s", url, leak)
			}
		}
	}
}

func ampOrQ(path string) string {
	if strings.Contains(path, "?") {
		return "&"
	}
	return "?"
}

// The live region must be outside every fragment that could replace it. A live
// region inside its own swap target is announced exactly never, which is the
// most expensive kind of accessibility bug: it looks correct in the markup and
// is silent in use.
func TestTheAnnouncerSurvivesEveryFragmentSwap(t *testing.T) {
	h := dashboard(t)

	page := get(t, h, "/")
	if !strings.Contains(page, `id="a11y-status"`) {
		t.Fatal("the page has no #a11y-status live region")
	}

	// Every swap target's fragment must not contain the announcer, or swapping
	// it would replace the node the screen reader is watching.
	for _, fragment := range []string{
		"/fragments/leaderboard",
		"/fragments/verdict",
		"/fragments/signals",
		"/fragments/service/pan",
	} {
		body := get(t, h, fragment)
		if strings.Contains(body, `id="a11y-status"`) {
			t.Errorf("%s contains the announcer, so swapping it replaces the live region", fragment)
		}
		if strings.Contains(body, `aria-live=`) {
			t.Errorf("%s declares its own aria-live region inside a swap target; it would be replaced rather than updated", fragment)
		}
	}
}

// Icons are glyphs in the shipped set, and a glyph is announced as whatever
// character it happens to be — "◐" is not "partial outage". The icon template
// hides them, which is correct, but then anything an icon is the only content of
// must carry a name some other way.
func TestNoControlIsNamedOnlyByAGlyph(t *testing.T) {
	h := dashboard(t)

	for _, path := range []string{"/", "/service/pan"} {
		problems := auditPath(t, h, path)
		for _, p := range problems {
			if p.Rule == "button-name" || p.Rule == "link-name" {
				t.Errorf("%s: %s", path, p)
			}
		}
	}
}
